# User Management Service

A complete user management system with authentication, authorization, and role-based access control.

## Features

- User registration and authentication
- JWT-based authentication
- Role-based access control (Admin/User roles)
- Password hashing with bcrypt
- RESTful API endpoints
- In-memory storage (easily extensible to database)

## Configuration

Add the following to your `config.yaml`:

```yaml
user_management:
  enabled: true
  http_port: "8080"
  jwt_secret: "change-this-secret-in-production"
  jwt_expiration_hours: 24
  default_admin_username: "admin"
  default_admin_email: "admin@example.com"
  default_admin_password: "admin123"
```

Or use environment variables:

```bash
export USER_MANAGEMENT_ENABLED=true
export USER_MANAGEMENT_HTTP_PORT=8080
export USER_MANAGEMENT_JWT_SECRET=your-secret-key
export USER_MANAGEMENT_JWT_EXPIRATION_HOURS=24
export USER_MANAGEMENT_DEFAULT_ADMIN_USERNAME=admin
export USER_MANAGEMENT_DEFAULT_ADMIN_EMAIL=admin@example.com
export USER_MANAGEMENT_DEFAULT_ADMIN_PASSWORD=admin123
```

## API Endpoints

### Public Endpoints

#### Register User
```bash
POST /api/users/register
Content-Type: application/json

{
  "username": "john",
  "email": "john@example.com",
  "password": "password123",
  "role": "user"
}
```

#### Login
```bash
POST /api/users/login
Content-Type: application/json

{
  "username": "john",
  "password": "password123"
}

Response:
{
  "token": "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...",
  "user": {
    "id": "uuid",
    "username": "john",
    "email": "john@example.com",
    "role": "user",
    "created_at": "2025-10-28T...",
    "updated_at": "2025-10-28T..."
  }
}
```

### Protected Endpoints

All protected endpoints require the `Authorization` header:

```
Authorization: Bearer <your-jwt-token>
```

#### List All Users (Admin Only)
```bash
GET /api/users
Authorization: Bearer <token>
```

#### Get User by ID
```bash
GET /api/users/:id
Authorization: Bearer <token>
```

#### Update User
```bash
PUT /api/users/:id
Authorization: Bearer <token>
Content-Type: application/json

{
  "username": "newusername",
  "email": "newemail@example.com",
  "role": "admin"
}
```

Note: Non-admin users can only update their own information and cannot change their role.

#### Delete User (Admin Only)
```bash
DELETE /api/users/:id
Authorization: Bearer <token>
```

#### Change Password
```bash
POST /api/users/change-password
Authorization: Bearer <token>
Content-Type: application/json

{
  "old_password": "oldpass123",
  "new_password": "newpass456"
}
```

## Usage Example

### 1. Login as default admin
```bash
curl -X POST http://localhost:8080/api/users/login \
  -H "Content-Type: application/json" \
  -d '{
    "username": "admin",
    "password": "admin123"
  }'
```

### 2. Create a new user
```bash
curl -X POST http://localhost:8080/api/users/register \
  -H "Content-Type: application/json" \
  -d '{
    "username": "newuser",
    "email": "newuser@example.com",
    "password": "password123",
    "role": "user"
  }'
```

### 3. List all users (admin only)
```bash
curl -X GET http://localhost:8080/api/users \
  -H "Authorization: Bearer <your-admin-token>"
```

### 4. Update user information
```bash
curl -X PUT http://localhost:8080/api/users/<user-id> \
  -H "Authorization: Bearer <your-token>" \
  -H "Content-Type: application/json" \
  -d '{
    "email": "updated@example.com"
  }'
```

### 5. Change password
```bash
curl -X POST http://localhost:8080/api/users/change-password \
  -H "Authorization: Bearer <your-token>" \
  -H "Content-Type: application/json" \
  -d '{
    "old_password": "oldpass123",
    "new_password": "newpass456"
  }'
```

## Security Best Practices

1. **Change Default Admin Password**: Always change the default admin credentials in production
2. **Use Strong JWT Secret**: Set a strong, random JWT secret key
3. **Use HTTPS**: Always use HTTPS in production to protect tokens and passwords in transit
4. **Secure Password Policy**: Enforce strong password requirements in your application
5. **Token Expiration**: Configure appropriate token expiration times based on your security needs

## Architecture

- **Models**: User data structures and request/response types
- **Storage**: In-memory storage with interface for easy database integration
- **Service**: Business logic for user management operations
- **Handlers**: HTTP request handlers for API endpoints
- **Middleware**: Authentication and authorization middleware
- **JWT**: Token generation and validation
- **Password**: Secure password hashing with bcrypt

## Future Enhancements

- Database persistence (PostgreSQL, MySQL, MongoDB)
- Email verification
- Password reset functionality
- Rate limiting
- Account lockout after failed login attempts
- OAuth2 integration
- Refresh tokens
- User sessions management
